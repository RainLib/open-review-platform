package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	minimumInvitationLifetime = 1
	maximumInvitationLifetime = 30 * 24
)

func invitationToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate invitation token: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func invitationTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func validInvitationInput(input domain.WorkspaceInvitationInput) bool {
	input.Subject = strings.TrimSpace(input.Subject)
	input.Role = strings.TrimSpace(input.Role)
	return input.Subject != "" && len(input.Subject) <= 240 &&
		!strings.ContainsAny(input.Subject, "\r\n\x00") &&
		domain.ValidInviteRole(input.Role) &&
		input.ExpiresInHours >= minimumInvitationLifetime &&
		input.ExpiresInHours <= maximumInvitationLifetime
}

func (s *PostgresStore) CreateWorkspaceInvitation(ctx context.Context, actor, tenantSlug string, input domain.WorkspaceInvitationInput) (domain.WorkspaceInvitationCreation, error) {
	input.Subject = strings.TrimSpace(input.Subject)
	input.Role = strings.TrimSpace(input.Role)
	if !validInvitationInput(input) {
		return domain.WorkspaceInvitationCreation{}, ErrInvalidInvitation
	}
	token, err := invitationToken()
	if err != nil {
		return domain.WorkspaceInvitationCreation{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.WorkspaceInvitationCreation{}, fmt.Errorf("begin workspace invitation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.WorkspaceInvitationCreation{}, err
	}
	if role != "owner" {
		return domain.WorkspaceInvitationCreation{}, ErrForbidden
	}
	var memberExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE tenant_id=$1 AND subject=$2)`, tenantID, input.Subject).Scan(&memberExists); err != nil {
		return domain.WorkspaceInvitationCreation{}, fmt.Errorf("check invited membership: %w", err)
	}
	if memberExists {
		return domain.WorkspaceInvitationCreation{}, ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE workspace_invitations SET status='expired' WHERE tenant_id=$1 AND subject=$2 AND status='pending' AND expires_at <= now()`, tenantID, input.Subject); err != nil {
		return domain.WorkspaceInvitationCreation{}, fmt.Errorf("expire previous workspace invitation: %w", err)
	}
	invitation := domain.WorkspaceInvitation{}
	err = tx.QueryRow(ctx, `
		INSERT INTO workspace_invitations (tenant_id,subject,role,token_sha256,created_by,expires_at)
		VALUES ($1,$2,$3,$4,$5,now()+($6 * interval '1 hour'))
		RETURNING id,tenant_id,subject,role,status,created_by,created_at,expires_at,accepted_at,accepted_by,revoked_at,revoked_by`,
		tenantID, input.Subject, input.Role, invitationTokenHash(token), actor, input.ExpiresInHours).Scan(
		&invitation.ID, &invitation.TenantID, &invitation.Subject, &invitation.Role,
		&invitation.Status, &invitation.CreatedBy, &invitation.CreatedAt, &invitation.ExpiresAt,
		&invitation.AcceptedAt, &invitation.AcceptedBy, &invitation.RevokedAt, &invitation.RevokedBy,
	)
	if isUniqueViolation(err) {
		return domain.WorkspaceInvitationCreation{}, ErrConflict
	}
	if err != nil {
		return domain.WorkspaceInvitationCreation{}, fmt.Errorf("create workspace invitation: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'workspace_invitation.created',$3,jsonb_build_object('subject',$4::text,'role',$5::text,'expires_at',$6::timestamptz))`, tenantID, actor, invitation.ID.String(), invitation.Subject, invitation.Role, invitation.ExpiresAt); err != nil {
		return domain.WorkspaceInvitationCreation{}, fmt.Errorf("audit workspace invitation creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.WorkspaceInvitationCreation{}, fmt.Errorf("commit workspace invitation: %w", err)
	}
	return domain.WorkspaceInvitationCreation{Invitation: invitation, Token: token}, nil
}

func (s *PostgresStore) ListWorkspaceInvitations(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.WorkspaceInvitation, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalidInvitation
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	if role != "owner" && role != "admin" {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id,tenant_id,subject,role,
		       CASE WHEN status='pending' AND expires_at <= now() THEN 'expired' ELSE status END,
		       created_by,created_at,expires_at,accepted_at,accepted_by,revoked_at,revoked_by
		FROM workspace_invitations WHERE tenant_id=$1
		ORDER BY created_at DESC,id DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list workspace invitations: %w", err)
	}
	defer rows.Close()
	items := make([]domain.WorkspaceInvitation, 0)
	for rows.Next() {
		var invitation domain.WorkspaceInvitation
		if err := rows.Scan(&invitation.ID, &invitation.TenantID, &invitation.Subject, &invitation.Role, &invitation.Status, &invitation.CreatedBy, &invitation.CreatedAt, &invitation.ExpiresAt, &invitation.AcceptedAt, &invitation.AcceptedBy, &invitation.RevokedAt, &invitation.RevokedBy); err != nil {
			return nil, fmt.Errorf("scan workspace invitation: %w", err)
		}
		items = append(items, invitation)
	}
	return items, rows.Err()
}

func (s *PostgresStore) AcceptWorkspaceInvitation(ctx context.Context, actor, token string) (domain.Membership, error) {
	actor = strings.TrimSpace(actor)
	token = strings.TrimSpace(token)
	if actor == "" || len(token) != 64 {
		return domain.Membership{}, ErrInvalidInvitation
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Membership{}, fmt.Errorf("begin workspace invitation acceptance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var invitation domain.WorkspaceInvitation
	err = tx.QueryRow(ctx, `
		SELECT id,tenant_id,subject,role,status,created_by,created_at,expires_at,accepted_at,accepted_by,revoked_at,revoked_by
		FROM workspace_invitations WHERE token_sha256=$1 FOR UPDATE`, invitationTokenHash(token)).Scan(
		&invitation.ID, &invitation.TenantID, &invitation.Subject, &invitation.Role, &invitation.Status,
		&invitation.CreatedBy, &invitation.CreatedAt, &invitation.ExpiresAt, &invitation.AcceptedAt,
		&invitation.AcceptedBy, &invitation.RevokedAt, &invitation.RevokedBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, ErrNotFound
	}
	if err != nil {
		return domain.Membership{}, fmt.Errorf("load workspace invitation: %w", err)
	}
	if invitation.Status != "pending" || invitation.ExpiresAt.Before(time.Now().UTC()) || invitation.Subject != actor {
		// Expiry is projected at read time and materialized before a subsequent
		// invite for the same subject. Do not write here: this rejection rolls
		// back its transaction and must not look like a durable state transition.
		return domain.Membership{}, ErrNotFound
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE tenant_id=$1 AND subject=$2)`, invitation.TenantID, actor).Scan(&exists); err != nil {
		return domain.Membership{}, fmt.Errorf("check accepted membership: %w", err)
	}
	if exists {
		return domain.Membership{}, ErrConflict
	}
	membership := domain.Membership{TenantID: invitation.TenantID, Subject: actor, Role: invitation.Role, Active: true}
	if _, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,$2,$3)`, membership.TenantID, membership.Subject, membership.Role); err != nil {
		return domain.Membership{}, fmt.Errorf("create invited membership: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE workspace_invitations SET status='accepted',accepted_at=now(),accepted_by=$2 WHERE id=$1`, invitation.ID, actor); err != nil {
		return domain.Membership{}, fmt.Errorf("accept workspace invitation: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'workspace_invitation.accepted',$3,jsonb_build_object('role',$4::text))`, invitation.TenantID, actor, invitation.ID.String(), invitation.Role); err != nil {
		return domain.Membership{}, fmt.Errorf("audit workspace invitation acceptance: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Membership{}, fmt.Errorf("commit workspace invitation acceptance: %w", err)
	}
	return membership, nil
}

func (s *PostgresStore) RevokeWorkspaceInvitation(ctx context.Context, actor, tenantSlug string, invitationID uuid.UUID) (domain.WorkspaceInvitation, error) {
	if invitationID == uuid.Nil {
		return domain.WorkspaceInvitation{}, ErrInvalidInvitation
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.WorkspaceInvitation{}, fmt.Errorf("begin workspace invitation revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.WorkspaceInvitation{}, err
	}
	if role != "owner" {
		return domain.WorkspaceInvitation{}, ErrForbidden
	}
	var invitation domain.WorkspaceInvitation
	err = tx.QueryRow(ctx, `
		UPDATE workspace_invitations SET status='revoked',revoked_at=now(),revoked_by=$3
		WHERE id=$1 AND tenant_id=$2 AND status='pending' AND expires_at > now()
		RETURNING id,tenant_id,subject,role,status,created_by,created_at,expires_at,accepted_at,accepted_by,revoked_at,revoked_by`, invitationID, tenantID, actor).Scan(
		&invitation.ID, &invitation.TenantID, &invitation.Subject, &invitation.Role, &invitation.Status,
		&invitation.CreatedBy, &invitation.CreatedAt, &invitation.ExpiresAt, &invitation.AcceptedAt,
		&invitation.AcceptedBy, &invitation.RevokedAt, &invitation.RevokedBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WorkspaceInvitation{}, ErrNotFound
	}
	if err != nil {
		return domain.WorkspaceInvitation{}, fmt.Errorf("revoke workspace invitation: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target) VALUES ($1,$2,'workspace_invitation.revoked',$3)`, tenantID, actor, invitation.ID.String()); err != nil {
		return domain.WorkspaceInvitation{}, fmt.Errorf("audit workspace invitation revocation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.WorkspaceInvitation{}, fmt.Errorf("commit workspace invitation revocation: %w", err)
	}
	return invitation, nil
}
