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

func (s *PostgresStore) ListIssueFormatTemplates(ctx context.Context, actor, tenantSlug string) ([]domain.IssueFormatTemplate, error) {
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT template.id, version.name, version.description, version.revision,
		       version.content, version.content_sha256,
		       template.created_by, template.created_at,
		       version.created_by, version.created_at
		FROM issue_format_templates template
		JOIN LATERAL (
			SELECT name, description, revision, content, content_sha256, created_by, created_at
			FROM issue_format_template_versions
			WHERE template_id=template.id
			ORDER BY revision DESC LIMIT 1
		) version ON TRUE
		WHERE template.tenant_id=$1 AND template.active=TRUE
		ORDER BY lower(version.name), template.id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list issue format templates: %w", err)
	}
	defer rows.Close()
	templates := make([]domain.IssueFormatTemplate, 0)
	for rows.Next() {
		var item domain.IssueFormatTemplate
		var content []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Revision,
			&content, &item.ContentSHA256, &item.CreatedBy, &item.CreatedAt,
			&item.UpdatedBy, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan issue format template: %w", err)
		}
		item.Content = json.RawMessage(content)
		templates = append(templates, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate issue format templates: %w", err)
	}
	return templates, nil
}

func (s *PostgresStore) CreateIssueFormatTemplate(ctx context.Context, actor, tenantSlug string, input domain.IssueFormatTemplateInput) (domain.IssueFormatTemplate, error) {
	normalized, hash, err := domain.NormalizeIssueFormatTemplateInput(input, false)
	if err != nil {
		return domain.IssueFormatTemplate{}, ErrInvalidIssueFormatTemplate
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.IssueFormatTemplate{}, fmt.Errorf("begin issue format template create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.IssueFormatTemplate{}, err
	}
	if !canManageIssueFormatTemplates(role) {
		return domain.IssueFormatTemplate{}, ErrForbidden
	}
	item := domain.IssueFormatTemplate{
		ID: uuid.New(), Name: normalized.Name, Description: normalized.Description,
		Revision: 1, Content: normalized.Content, ContentSHA256: hash,
		CreatedBy: actor, UpdatedBy: actor,
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO issue_format_templates (id,tenant_id,name_key,created_by)
		VALUES ($1,$2,$3,$4)
		RETURNING created_at,updated_at`, item.ID, tenantID, domain.IssueFormatTemplateNameKey(item.Name), actor).Scan(&item.CreatedAt, &item.UpdatedAt); isUniqueViolation(err) {
		return domain.IssueFormatTemplate{}, ErrConflict
	} else if err != nil {
		return domain.IssueFormatTemplate{}, fmt.Errorf("create issue format template: %w", err)
	}
	if err := insertIssueFormatTemplateVersion(ctx, tx, item.ID, item.Revision, normalized, hash, actor); err != nil {
		return domain.IssueFormatTemplate{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'issue_format_template.created',$3,
		        jsonb_build_object('revision',1,'content_sha256',$4::text,'name',$5::text))`, tenantID, actor, item.ID.String(), hash, item.Name); err != nil {
		return domain.IssueFormatTemplate{}, fmt.Errorf("audit issue format template create: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.IssueFormatTemplate{}, fmt.Errorf("commit issue format template create: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) UpdateIssueFormatTemplate(ctx context.Context, actor, tenantSlug string, templateID uuid.UUID, input domain.IssueFormatTemplateInput) (domain.IssueFormatTemplate, error) {
	if templateID == uuid.Nil {
		return domain.IssueFormatTemplate{}, ErrInvalidIssueFormatTemplate
	}
	normalized, hash, err := domain.NormalizeIssueFormatTemplateInput(input, true)
	if err != nil {
		return domain.IssueFormatTemplate{}, ErrInvalidIssueFormatTemplate
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.IssueFormatTemplate{}, fmt.Errorf("begin issue format template update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.IssueFormatTemplate{}, err
	}
	if !canManageIssueFormatTemplates(role) {
		return domain.IssueFormatTemplate{}, ErrForbidden
	}
	item, err := lockIssueFormatTemplate(ctx, tx, tenantID, templateID)
	if err != nil {
		return domain.IssueFormatTemplate{}, err
	}
	if item.Revision != normalized.ExpectedRevision {
		return domain.IssueFormatTemplate{}, ErrRevisionConflict
	}
	item.Name, item.Description = normalized.Name, normalized.Description
	item.Revision++
	item.Content, item.ContentSHA256 = normalized.Content, hash
	item.UpdatedBy = actor
	if _, err := tx.Exec(ctx, `UPDATE issue_format_templates SET name_key=$2,updated_at=now() WHERE id=$1`, templateID, domain.IssueFormatTemplateNameKey(item.Name)); isUniqueViolation(err) {
		return domain.IssueFormatTemplate{}, ErrConflict
	} else if err != nil {
		return domain.IssueFormatTemplate{}, fmt.Errorf("update issue format template identity: %w", err)
	}
	if err := insertIssueFormatTemplateVersion(ctx, tx, templateID, item.Revision, normalized, hash, actor); err != nil {
		return domain.IssueFormatTemplate{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT updated_at FROM issue_format_templates WHERE id=$1`, templateID).Scan(&item.UpdatedAt); err != nil {
		return domain.IssueFormatTemplate{}, fmt.Errorf("read issue format template update time: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'issue_format_template.updated',$3,
		        jsonb_build_object('revision',$4::integer,'content_sha256',$5::text,'name',$6::text))`, tenantID, actor, templateID.String(), item.Revision, hash, item.Name); err != nil {
		return domain.IssueFormatTemplate{}, fmt.Errorf("audit issue format template update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.IssueFormatTemplate{}, fmt.Errorf("commit issue format template update: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) ArchiveIssueFormatTemplate(ctx context.Context, actor, tenantSlug string, templateID uuid.UUID, expectedRevision int) error {
	if templateID == uuid.Nil || expectedRevision < 1 {
		return ErrInvalidIssueFormatTemplate
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin issue format template archive: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return err
	}
	if !canManageIssueFormatTemplates(role) {
		return ErrForbidden
	}
	item, err := lockIssueFormatTemplate(ctx, tx, tenantID, templateID)
	if err != nil {
		return err
	}
	if item.Revision != expectedRevision {
		return ErrRevisionConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE issue_format_templates SET active=FALSE,updated_at=now() WHERE id=$1`, templateID); err != nil {
		return fmt.Errorf("archive issue format template: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'issue_format_template.archived',$3,
		        jsonb_build_object('revision',$4::integer,'content_sha256',$5::text,'name',$6::text))`, tenantID, actor, templateID.String(), item.Revision, item.ContentSHA256, item.Name); err != nil {
		return fmt.Errorf("audit issue format template archive: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit issue format template archive: %w", err)
	}
	return nil
}

func lockIssueFormatTemplate(ctx context.Context, tx pgx.Tx, tenantID, templateID uuid.UUID) (domain.IssueFormatTemplate, error) {
	var item domain.IssueFormatTemplate
	var content []byte
	err := tx.QueryRow(ctx, `
		SELECT template.id, version.name, version.description, version.revision,
		       version.content, version.content_sha256,
		       template.created_by, template.created_at,
		       version.created_by, version.created_at
		FROM issue_format_templates template
		JOIN LATERAL (
			SELECT name, description, revision, content, content_sha256, created_by, created_at
			FROM issue_format_template_versions
			WHERE template_id=template.id ORDER BY revision DESC LIMIT 1
		) version ON TRUE
		WHERE template.id=$1 AND template.tenant_id=$2 AND template.active=TRUE
		FOR UPDATE OF template`, templateID, tenantID).Scan(
		&item.ID, &item.Name, &item.Description, &item.Revision,
		&content, &item.ContentSHA256, &item.CreatedBy, &item.CreatedAt,
		&item.UpdatedBy, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.IssueFormatTemplate{}, ErrNotFound
	}
	if err != nil {
		return domain.IssueFormatTemplate{}, fmt.Errorf("lock issue format template: %w", err)
	}
	item.Content = json.RawMessage(content)
	return item, nil
}

func insertIssueFormatTemplateVersion(ctx context.Context, tx pgx.Tx, templateID uuid.UUID, revision int, input domain.IssueFormatTemplateInput, hash, actor string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO issue_format_template_versions
		       (template_id,revision,name,description,content,content_sha256,created_by)
		VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7)`, templateID, revision, input.Name, input.Description, input.Content, hash, actor)
	if err != nil {
		return fmt.Errorf("version issue format template: %w", err)
	}
	return nil
}

func canManageIssueFormatTemplates(role string) bool {
	return role == "owner" || role == "admin" || role == "rule_admin"
}
