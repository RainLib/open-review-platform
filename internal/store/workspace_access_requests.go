package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var accessRequestSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

const accessRequestColumns = `id, tenant_id, subject, note, status, revision, created_at, decided_at, decided_by`

func validAccessRequestInput(actor, slug, note string) bool {
	return actor != "" && len(actor) <= 240 && !strings.ContainsAny(actor, "\r\n\x00") &&
		accessRequestSlugPattern.MatchString(slug) && len(note) <= 1000 &&
		!strings.ContainsAny(note, "\x00")
}

// RequestWorkspaceAccess deliberately returns the same result for an unknown
// slug, existing member, duplicate, or throttled request. Only an authenticated
// owner can subsequently see a durable request for their own workspace.
func (s *PostgresStore) RequestWorkspaceAccess(ctx context.Context, actor, tenantSlug, note string) error {
	actor, tenantSlug, note = strings.TrimSpace(actor), strings.TrimSpace(tenantSlug), strings.TrimSpace(note)
	if !validAccessRequestInput(actor, tenantSlug, note) {
		return ErrInvalidAccessRequest
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin workspace access request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize a requester's cross-workspace throttle, including concurrent
	// requests for different slugs. No slug or existence result is returned.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, actor); err != nil {
		return fmt.Errorf("lock workspace access request actor: %w", err)
	}
	var tenantID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM tenants WHERE slug=$1`, tenantSlug).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve workspace access request: %w", err)
	}
	var alreadyMember, pending, recent, throttled bool
	err = tx.QueryRow(ctx, `
		SELECT
		  EXISTS(SELECT 1 FROM memberships WHERE tenant_id=$1 AND subject=$2),
		  EXISTS(SELECT 1 FROM workspace_access_requests WHERE tenant_id=$1 AND subject=$2 AND status='pending'),
		  EXISTS(SELECT 1 FROM workspace_access_requests WHERE tenant_id=$1 AND subject=$2 AND created_at>now()-interval '24 hours'),
		  (SELECT count(*)>=5 FROM workspace_access_requests WHERE subject=$2 AND created_at>now()-interval '24 hours')`, tenantID, actor).Scan(&alreadyMember, &pending, &recent, &throttled)
	if err != nil {
		return fmt.Errorf("check workspace access request limits: %w", err)
	}
	if alreadyMember || pending || recent || throttled {
		return nil
	}
	var requestID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO workspace_access_requests(tenant_id,subject,note)
		VALUES($1,$2,$3)
		ON CONFLICT (tenant_id,subject) WHERE status='pending' DO NOTHING
		RETURNING id`, tenantID, actor, note).Scan(&requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create workspace access request: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target) VALUES($1,$2,'workspace_access.requested',$3)`, tenantID, actor, requestID.String()); err != nil {
		return fmt.Errorf("audit workspace access request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit workspace access request: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListWorkspaceAccessRequests(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.WorkspaceAccessRequest, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalidAccessRequest
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	if role != "owner" && role != "admin" {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT `+accessRequestColumns+` FROM workspace_access_requests WHERE tenant_id=$1 AND status='pending' ORDER BY created_at DESC,id DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list workspace access requests: %w", err)
	}
	defer rows.Close()
	items := make([]domain.WorkspaceAccessRequest, 0)
	for rows.Next() {
		item, scanErr := scanWorkspaceAccessRequest(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan workspace access request: %w", scanErr)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) DecideWorkspaceAccessRequest(ctx context.Context, actor, tenantSlug string, requestID uuid.UUID, expectedRevision int, decision string) (domain.WorkspaceAccessRequest, error) {
	if requestID == uuid.Nil || expectedRevision < 1 || (decision != "approve" && decision != "reject") {
		return domain.WorkspaceAccessRequest{}, ErrInvalidAccessRequest
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.WorkspaceAccessRequest{}, fmt.Errorf("begin workspace access decision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.WorkspaceAccessRequest{}, err
	}
	if role != "owner" {
		return domain.WorkspaceAccessRequest{}, ErrForbidden
	}
	item, err := scanWorkspaceAccessRequest(tx.QueryRow(ctx, `SELECT `+accessRequestColumns+` FROM workspace_access_requests WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, requestID, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WorkspaceAccessRequest{}, ErrNotFound
	}
	if err != nil {
		return domain.WorkspaceAccessRequest{}, fmt.Errorf("load workspace access request: %w", err)
	}
	if item.Status != "pending" || item.Revision != expectedRevision {
		return domain.WorkspaceAccessRequest{}, ErrRevisionConflict
	}
	if decision == "approve" {
		// A request grants only viewer access. Existing memberships, including
		// deactivated ones, require the explicit membership management flow.
		result, insertErr := tx.Exec(ctx, `INSERT INTO memberships(tenant_id,subject,role) VALUES($1,$2,'viewer') ON CONFLICT (tenant_id,subject) DO NOTHING`, tenantID, item.Subject)
		if insertErr != nil {
			return domain.WorkspaceAccessRequest{}, fmt.Errorf("grant requested workspace access: %w", insertErr)
		}
		if result.RowsAffected() != 1 {
			return domain.WorkspaceAccessRequest{}, ErrConflict
		}
	}
	status := "rejected"
	grantedRole := ""
	if decision == "approve" {
		status = "approved"
		grantedRole = "viewer"
	}
	item, err = scanWorkspaceAccessRequest(tx.QueryRow(ctx, `UPDATE workspace_access_requests SET status=$3,revision=revision+1,decided_at=now(),decided_by=$4 WHERE id=$1 AND tenant_id=$2 RETURNING `+accessRequestColumns, requestID, tenantID, status, actor))
	if err != nil {
		return domain.WorkspaceAccessRequest{}, fmt.Errorf("record workspace access decision: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'workspace_access.decided',$3,jsonb_build_object('decision',$4::text,'subject',$5::text,'role',$6::text))`, tenantID, actor, item.ID.String(), decision, item.Subject, grantedRole); err != nil {
		return domain.WorkspaceAccessRequest{}, fmt.Errorf("audit workspace access decision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.WorkspaceAccessRequest{}, fmt.Errorf("commit workspace access decision: %w", err)
	}
	return item, nil
}

func scanWorkspaceAccessRequest(row rowScanner) (domain.WorkspaceAccessRequest, error) {
	var item domain.WorkspaceAccessRequest
	err := row.Scan(&item.ID, &item.TenantID, &item.Subject, &item.Note, &item.Status, &item.Revision, &item.CreatedAt, &item.DecidedAt, &item.DecidedBy)
	return item, err
}
