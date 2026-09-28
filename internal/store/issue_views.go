package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const issueViewColumns = `id,name,visibility,revision,definition,owner_subject,created_at,updated_at`

func (s *PostgresStore) ListIssueViews(ctx context.Context, actor, slug string) (domain.IssueSavedViewPage, error) {
	tenantID, role, err := s.authorizedTenant(ctx, actor, slug)
	if err != nil {
		return domain.IssueSavedViewPage{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+issueViewColumns+` FROM issue_saved_views WHERE tenant_id=$1 AND (visibility='workspace' OR owner_subject=$2) ORDER BY visibility,name,id LIMIT 201`, tenantID, actor)
	if err != nil {
		return domain.IssueSavedViewPage{}, fmt.Errorf("list saved issue views: %w", err)
	}
	defer rows.Close()
	page := domain.IssueSavedViewPage{Views: []domain.IssueSavedView{}, CanCreateWorkspace: canManageWorkspaceIssueViews(role)}
	for rows.Next() {
		view, err := scanIssueView(rows)
		if err != nil {
			return domain.IssueSavedViewPage{}, err
		}
		view.CanManage = canManageIssueView(actor, role, view)
		page.Views = append(page.Views, view)
	}
	if err := rows.Err(); err != nil {
		return domain.IssueSavedViewPage{}, fmt.Errorf("iterate saved issue views: %w", err)
	}
	if len(page.Views) > 200 {
		return domain.IssueSavedViewPage{}, ErrConflict // Never return a silently truncated directory.
	}
	return page, nil
}

func (s *PostgresStore) CreateIssueView(ctx context.Context, actor, slug string, input domain.IssueSavedViewInput) (domain.IssueSavedView, error) {
	return s.writeIssueView(ctx, actor, slug, uuid.Nil, input)
}

func (s *PostgresStore) UpdateIssueView(ctx context.Context, actor, slug string, id uuid.UUID, input domain.IssueSavedViewInput) (domain.IssueSavedView, error) {
	if id == uuid.Nil {
		return domain.IssueSavedView{}, ErrInvalidIssueFilter
	}
	return s.writeIssueView(ctx, actor, slug, id, input)
}

func (s *PostgresStore) writeIssueView(ctx context.Context, actor, slug string, id uuid.UUID, input domain.IssueSavedViewInput) (domain.IssueSavedView, error) {
	input.Name = strings.TrimSpace(input.Name)
	if !input.Valid() || (id == uuid.Nil && input.Revision != 0) || (id != uuid.Nil && input.Revision < 1) {
		return domain.IssueSavedView{}, ErrInvalidIssueFilter
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.IssueSavedView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, slug)
	if err != nil {
		return domain.IssueSavedView{}, err
	}
	// Serialize directory mutations to enforce per-owner/shared caps even when
	// simultaneous create and visibility-change requests race.
	if _, err := tx.Exec(ctx, `SELECT id FROM tenants WHERE id=$1 FOR UPDATE`, tenantID); err != nil {
		return domain.IssueSavedView{}, err
	}
	owner, action := actor, "issue_view.created"
	if id != uuid.Nil {
		existing, err := lockIssueView(ctx, tx, tenantID, id, actor)
		if err != nil {
			return domain.IssueSavedView{}, err
		}
		if !canManageIssueView(actor, role, existing) {
			return domain.IssueSavedView{}, ErrForbidden
		}
		if existing.Revision != input.Revision {
			return domain.IssueSavedView{}, ErrRevisionConflict
		}
		owner, action = existing.OwnerSubject, "issue_view.updated"
		if existing.Visibility != input.Visibility && input.Visibility == "personal" {
			owner = actor
		}
	}
	if input.Visibility == "workspace" && !canManageWorkspaceIssueViews(role) {
		return domain.IssueSavedView{}, ErrForbidden
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM issue_saved_views WHERE tenant_id=$1 AND visibility=$2 AND ($2='workspace' OR owner_subject=$3) AND id<>$4`, tenantID, input.Visibility, owner, id).Scan(&count); err != nil {
		return domain.IssueSavedView{}, err
	}
	if count >= 100 {
		return domain.IssueSavedView{}, ErrConflict
	}
	definition, err := json.Marshal(input.Definition)
	if err != nil {
		return domain.IssueSavedView{}, ErrInvalidIssueFilter
	}
	var result domain.IssueSavedView
	if id == uuid.Nil {
		result, err = scanIssueView(tx.QueryRow(ctx, `INSERT INTO issue_saved_views(tenant_id,owner_subject,visibility,name,definition) VALUES($1,$2,$3,$4,$5) RETURNING `+issueViewColumns, tenantID, owner, input.Visibility, input.Name, definition))
	} else {
		result, err = scanIssueView(tx.QueryRow(ctx, `UPDATE issue_saved_views SET owner_subject=$3,visibility=$4,name=$5,definition=$6,revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2 RETURNING `+issueViewColumns, tenantID, id, owner, input.Visibility, input.Name, definition))
	}
	if isUniqueViolation(err) {
		return domain.IssueSavedView{}, ErrConflict
	}
	if err != nil {
		return domain.IssueSavedView{}, fmt.Errorf("save issue view: %w", err)
	}
	if err := auditIssueView(ctx, tx, tenantID, actor, action, result); err != nil {
		return domain.IssueSavedView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.IssueSavedView{}, err
	}
	result.CanManage = true
	return result, nil
}

func (s *PostgresStore) DeleteIssueView(ctx context.Context, actor, slug string, id uuid.UUID, revision int) error {
	if id == uuid.Nil || revision < 1 {
		return ErrInvalidIssueFilter
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, slug)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM tenants WHERE id=$1 FOR UPDATE`, tenantID); err != nil {
		return err
	}
	view, err := lockIssueView(ctx, tx, tenantID, id, actor)
	if err != nil {
		return err
	}
	if !canManageIssueView(actor, role, view) {
		return ErrForbidden
	}
	if view.Revision != revision {
		return ErrRevisionConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM issue_saved_views WHERE id=$1 AND tenant_id=$2`, id, tenantID); err != nil {
		return err
	}
	if err := auditIssueView(ctx, tx, tenantID, actor, "issue_view.deleted", view); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockIssueView(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID, actor string) (domain.IssueSavedView, error) {
	view, err := scanIssueView(tx.QueryRow(ctx, `SELECT `+issueViewColumns+` FROM issue_saved_views WHERE tenant_id=$1 AND id=$2 AND (visibility='workspace' OR owner_subject=$3) FOR UPDATE`, tenantID, id, actor))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.IssueSavedView{}, ErrNotFound
	}
	return view, err
}

func scanIssueView(row rowScanner) (domain.IssueSavedView, error) {
	var view domain.IssueSavedView
	var definition []byte
	if err := row.Scan(&view.ID, &view.Name, &view.Visibility, &view.Revision, &definition, &view.OwnerSubject, &view.CreatedAt, &view.UpdatedAt); err != nil {
		return domain.IssueSavedView{}, err
	}
	if err := json.Unmarshal(definition, &view.Definition); err != nil {
		return domain.IssueSavedView{}, fmt.Errorf("decode issue view: %w", err)
	}
	return view, nil
}

func auditIssueView(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, actor, action string, view domain.IssueSavedView) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,$3,$4,jsonb_build_object('revision',$5::integer,'visibility',$6::text,'name',$7::text))`, tenantID, actor, action, view.ID.String(), view.Revision, view.Visibility, view.Name)
	return err
}

func canManageWorkspaceIssueViews(role string) bool { return role == "owner" || role == "admin" }
func canManageIssueView(actor, role string, view domain.IssueSavedView) bool {
	return (view.Visibility == "personal" && view.OwnerSubject == actor) || (view.Visibility == "workspace" && canManageWorkspaceIssueViews(role))
}
