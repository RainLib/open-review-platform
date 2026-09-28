package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const apiKeyTokenPrefix = "orp_live_"

func (s *PostgresStore) CreateAPIKey(ctx context.Context, actor, tenantSlug string, input domain.APIKeyInput) (domain.APIKeyCreation, error) {
	input, valid := domain.NormalizeAPIKeyInput(input, time.Now())
	if !valid {
		return domain.APIKeyCreation{}, ErrInvalidAPIKey
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.APIKeyCreation{}, fmt.Errorf("begin API key creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.APIKeyCreation{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.APIKeyCreation{}, ErrForbidden
	}
	secret, prefix, hash, err := generateAPIKeySecret()
	if err != nil {
		return domain.APIKeyCreation{}, err
	}
	item := domain.APIKey{
		ID: uuid.New(), Name: input.Name, Prefix: prefix, Scopes: input.Scopes,
		Repositories: input.Repositories, CreatedBy: actor, ExpiresAt: input.ExpiresAt,
	}
	item.CallerSubject = "api-key:" + item.ID.String()
	err = tx.QueryRow(ctx, `
		INSERT INTO api_keys (
			id, tenant_id, name, key_prefix, secret_hash, caller_subject,
			scopes, repositories, created_by, expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING created_at`, item.ID, tenantID, input.Name, prefix, hash[:], item.CallerSubject, input.Scopes, input.Repositories, actor, input.ExpiresAt).
		Scan(&item.CreatedAt)
	if err != nil {
		return domain.APIKeyCreation{}, fmt.Errorf("create API key: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1, $2, 'api_key.created', $3, jsonb_build_object(
			'name', $4::text, 'prefix', $5::text, 'scopes', $6::text[],
			'repositories', $7::text[], 'expires_at', $8::timestamptz
		))`, tenantID, actor, item.ID.String(), item.Name, item.Prefix, item.Scopes, item.Repositories, item.ExpiresAt); err != nil {
		return domain.APIKeyCreation{}, fmt.Errorf("audit API key creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.APIKeyCreation{}, fmt.Errorf("commit API key creation: %w", err)
	}
	return domain.APIKeyCreation{APIKey: item, Secret: secret}, nil
}

func (s *PostgresStore) ListAPIKeys(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.APIKey, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalidAPIKey
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	if role != "owner" && role != "admin" {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, key_prefix, caller_subject, scopes, repositories,
		       created_by, created_at, expires_at, last_used_at, revoked_at, revoked_by
		FROM api_keys
		WHERE tenant_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list API keys: %w", err)
	}
	defer rows.Close()
	items := make([]domain.APIKey, 0)
	for rows.Next() {
		item, err := scanAPIKey(rows)
		if err != nil {
			return nil, fmt.Errorf("scan API key: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) RevokeAPIKey(ctx context.Context, actor, tenantSlug string, keyID uuid.UUID) (domain.APIKey, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.APIKey{}, fmt.Errorf("begin API key revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.APIKey{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.APIKey{}, ErrForbidden
	}
	item, err := scanAPIKey(tx.QueryRow(ctx, `
		UPDATE api_keys
		SET revoked_at = COALESCE(revoked_at, now()),
		    revoked_by = CASE WHEN revoked_at IS NULL THEN $3 ELSE revoked_by END
		WHERE id = $1 AND tenant_id = $2
		RETURNING id, name, key_prefix, caller_subject, scopes, repositories,
		          created_by, created_at, expires_at, last_used_at, revoked_at, revoked_by`, keyID, tenantID, actor))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.APIKey{}, ErrNotFound
	}
	if err != nil {
		return domain.APIKey{}, fmt.Errorf("revoke API key: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1, $2, 'api_key.revoked', $3, jsonb_build_object('prefix', $4::text))`,
		tenantID, actor, item.ID.String(), item.Prefix); err != nil {
		return domain.APIKey{}, fmt.Errorf("audit API key revocation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.APIKey{}, fmt.Errorf("commit API key revocation: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) AuthenticateAPIKey(ctx context.Context, secret string) (domain.APIKeyPrincipal, error) {
	secret = strings.TrimSpace(secret)
	if !strings.HasPrefix(secret, apiKeyTokenPrefix) || len(secret) < len(apiKeyTokenPrefix)+40 || len(secret) > 100 {
		return domain.APIKeyPrincipal{}, ErrInvalidAPIKeyCredential
	}
	hash := sha256.Sum256([]byte(secret))
	var principal domain.APIKeyPrincipal
	err := s.pool.QueryRow(ctx, `
		UPDATE api_keys key
		SET last_used_at = now()
		FROM tenants tenant
		WHERE key.tenant_id = tenant.id
		  AND key.secret_hash = $1
		  AND key.revoked_at IS NULL
		  AND (key.expires_at IS NULL OR key.expires_at > now())
		RETURNING key.id, key.tenant_id, tenant.slug, key.caller_subject,
		          key.scopes, key.repositories`, hash[:]).Scan(
		&principal.KeyID, &principal.TenantID, &principal.TenantSlug,
		&principal.Subject, &principal.Scopes, &principal.Repositories,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.APIKeyPrincipal{}, ErrInvalidAPIKeyCredential
	}
	if err != nil {
		return domain.APIKeyPrincipal{}, fmt.Errorf("authenticate API key: %w", err)
	}
	return principal, nil
}

func generateAPIKeySecret() (secret string, prefix string, hash [32]byte, err error) {
	random := make([]byte, 32)
	if _, err = rand.Read(random); err != nil {
		return "", "", hash, fmt.Errorf("generate API key secret: %w", err)
	}
	secret = apiKeyTokenPrefix + base64.RawURLEncoding.EncodeToString(random)
	prefix = secret[:20]
	hash = sha256.Sum256([]byte(secret))
	return secret, prefix, hash, nil
}

type apiKeyScanner interface {
	Scan(dest ...any) error
}

func scanAPIKey(row apiKeyScanner) (domain.APIKey, error) {
	var item domain.APIKey
	err := row.Scan(
		&item.ID, &item.Name, &item.Prefix, &item.CallerSubject,
		&item.Scopes, &item.Repositories, &item.CreatedBy, &item.CreatedAt,
		&item.ExpiresAt, &item.LastUsedAt, &item.RevokedAt, &item.RevokedBy,
	)
	return item, err
}
